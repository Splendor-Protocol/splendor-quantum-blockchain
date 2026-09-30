package state

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// withRootConfig runs fn with the reducer's chunk size and worker count forced,
// then restores them.
func withRootConfig(t *testing.T, chunk, workers int, fn func()) {
	t.Helper()
	oldChunk, oldWorkers := rootChunkLeaves, rootWorkers
	rootChunkLeaves = chunk
	rootWorkers = func() int { return workers }
	defer func() { rootChunkLeaves, rootWorkers = oldChunk, oldWorkers }()
	fn()
}

// randIndex returns n leaves with random binary keys of varied length, keyed
// the way the state index is (key -> SHA3-256 of the value).
func randIndex(rng *rand.Rand, n int) map[string][32]byte {
	idx := make(map[string][32]byte, n)
	for len(idx) < n {
		k := make([]byte, 1+rng.Intn(40))
		rng.Read(k)
		var v [32]byte
		rng.Read(v[:])
		idx[string(k)] = v
	}
	return idx
}

func sortedKeys(idx map[string][32]byte) []string {
	keys := make([]string, 0, len(idx))
	for k := range idx {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mustApply(t *testing.T, tr *leafTree, delta []leafChange) *leafTree {
	t.Helper()
	out, err := tr.apply(delta)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustTree(t *testing.T, idx map[string][32]byte) *leafTree {
	t.Helper()
	tr, err := leafTreeFromIndex(idx)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// The tree shape written out by hand: leaves in key order, pairs hashed left
// to right, an odd last node carried up unchanged. This pins the construction
// both merkleRoot and the tree must reproduce.
func TestLeafTreeRootByHand(t *testing.T) {
	v := func(s string) [32]byte { return qcrypto.Hash32([]byte(s)) }
	idx := map[string][32]byte{
		"acc:e": v("5"), "acc:a": v("1"), "code:c": v("3"), "acc:b": v("2"), "store:d": v("4"),
	}
	d := func(k string) [32]byte { return leafDigest(k, idx[k]) }
	// Key order: acc:a acc:b acc:e code:c store:d.
	a, b, e, c, s := d("acc:a"), d("acc:b"), d("acc:e"), d("code:c"), d("store:d")

	cases := []struct {
		keys []string
		want [32]byte
	}{
		{nil, stateEmptyRoot},
		{[]string{"acc:a"}, a},
		{[]string{"acc:a", "acc:b"}, internalHash(a, b)},
		{[]string{"acc:a", "acc:b", "acc:e"}, internalHash(internalHash(a, b), e)},
		{[]string{"acc:a", "acc:b", "acc:e", "code:c", "store:d"},
			internalHash(internalHash(internalHash(a, b), internalHash(e, c)), s)},
	}
	for _, tc := range cases {
		sub := map[string][32]byte{}
		for _, k := range tc.keys {
			sub[k] = idx[k]
		}
		if got := merkleRoot(sub); got != tc.want {
			t.Fatalf("merkleRoot over %d leaves = %x, want %x", len(tc.keys), got[:8], tc.want[:8])
		}
		withRootConfig(t, 2, 3, func() {
			if got := mustTree(t, sub).root; got != tc.want {
				t.Fatalf("tree over %d leaves = %x, want %x", len(tc.keys), got[:8], tc.want[:8])
			}
		})
	}
}

// Chunked, parallel reduction must equal the sequential reference at every
// size, including each chunk boundary and its neighbours.
func TestLeafTreeRootMatchesReference(t *testing.T) {
	var sizes []int
	for n := 0; n <= 300; n++ {
		sizes = append(sizes, n)
	}
	sizes = append(sizes, 511, 512, 513, 1023, 1024, 1025, 1100)
	rng := rand.New(rand.NewSource(7))
	for _, n := range sizes {
		idx := randIndex(rng, n)
		want := merkleRoot(idx)
		for _, chunk := range []int{2, 4, 8, 4096} {
			for workers := 1; workers <= 4; workers++ {
				withRootConfig(t, chunk, workers, func() {
					if got := mustTree(t, idx).root; got != want {
						t.Fatalf("n=%d chunk=%d workers=%d: %x, want %x", n, chunk, workers, got[:8], want[:8])
					}
				})
			}
		}
	}
}

// A later write to the same key wins, as it does in the commit batch, and the
// change set comes out sorted by key with each leaf's digest.
func TestChangeSetLastWriteWinsInKeyOrder(t *testing.T) {
	h := func(s string) [32]byte { return qcrypto.Hash32([]byte(s)) }
	got := changeSet([]leafWrite{
		{key: "store:b", hash: h("old")},
		{key: "acc:z", hash: h("z")},
		{key: "store:b", hash: h("new")},
		{key: "acc:a", hash: h("a")},
	})
	want := []leafChange{
		{key: "acc:a", dig: leafDigest("acc:a", h("a"))},
		{key: "acc:z", dig: leafDigest("acc:z", h("z"))},
		{key: "store:b", dig: leafDigest("store:b", h("new"))},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d changes, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("change %d = %q %x, want %q %x", i, got[i].key, got[i].dig[:4], want[i].key, want[i].dig[:4])
		}
	}
	if len(changeSet(nil)) != 0 {
		t.Fatal("no writes must give an empty change set")
	}
}

// checkApplied verifies a tree produced by apply against the merged index:
// same keys in order, same root as the reference.
func checkApplied(t *testing.T, name string, got *leafTree, merged map[string][32]byte) {
	t.Helper()
	keys := sortedKeys(merged)
	if got.size() != len(keys) {
		t.Fatalf("%s: %d leaves, want %d", name, got.size(), len(keys))
	}
	for i, k := range keys {
		if got.key(i) != k {
			t.Fatalf("%s: key %d = %q, want %q", name, i, got.key(i), k)
		}
		if got.dig[i] != leafDigest(k, merged[k]) {
			t.Fatalf("%s: digest %d is stale", name, i)
		}
	}
	if want := merkleRoot(merged); got.root != want {
		t.Fatalf("%s: root %x, want %x", name, got.root[:8], want[:8])
	}
}

func TestLeafTreeApplyEdges(t *testing.T) {
	h := func(s string) [32]byte { return qcrypto.Hash32([]byte(s)) }
	base := map[string][32]byte{"b": h("b"), "d": h("d"), "f": h("f"), "h": h("h"), "j": h("j")}
	cases := []struct {
		name string
		base map[string][32]byte
		set  map[string][32]byte
	}{
		{"insert before the first key", base, map[string][32]byte{"a": h("a")}},
		{"insert after the last key", base, map[string][32]byte{"z": h("z")}},
		{"adjacent inserts", base, map[string][32]byte{"c": h("c"), "ca": h("ca"), "cb": h("cb")}},
		{"insert next to an update", base, map[string][32]byte{"e": h("e"), "f": h("f2")}},
		{"update every key", base, map[string][32]byte{"b": h("1"), "d": h("2"), "f": h("3"), "h": h("4"), "j": h("5")}},
		{"rewrite a key with its own value", base, map[string][32]byte{"d": h("d")}},
		{"inserts into an empty tree", map[string][32]byte{}, map[string][32]byte{"m": h("m"), "a": h("a")}},
		{"one insert into a one-leaf tree", map[string][32]byte{"k": h("k")}, map[string][32]byte{"a": h("a")}},
	}
	for _, tc := range cases {
		withRootConfig(t, 2, 2, func() {
			tr := mustTree(t, tc.base)
			baseRoot := tr.root
			var writes []leafWrite
			merged := map[string][32]byte{}
			for k, v := range tc.base {
				merged[k] = v
			}
			for k, v := range tc.set {
				writes = append(writes, leafWrite{key: k, hash: v})
				merged[k] = v
			}
			got := mustApply(t, tr, changeSet(writes))
			checkApplied(t, tc.name, got, merged)
			// The base tree is immutable: readers holding it see no change.
			checkApplied(t, tc.name+" (base after apply)", tr, tc.base)
			if tr.root != baseRoot {
				t.Fatalf("%s: apply moved the base root", tc.name)
			}
		})
	}
}

// Random change sets — inserts and updates at random positions — applied to
// random trees must always land on the reference root.
func TestLeafTreeApplyRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for iter := 0; iter < 400; iter++ {
		n := rng.Intn(600)
		chunk := []int{2, 4, 8, 4096}[rng.Intn(4)]
		workers := 1 + rng.Intn(4)
		withRootConfig(t, chunk, workers, func() {
			base := randIndex(rng, n)
			tr := mustTree(t, base)
			merged := map[string][32]byte{}
			for k, v := range base {
				merged[k] = v
			}
			var writes []leafWrite
			keys := sortedKeys(base)
			for c := rng.Intn(12); c > 0; c-- {
				var v [32]byte
				rng.Read(v[:])
				k := ""
				if len(keys) > 0 && rng.Intn(2) == 0 {
					k = keys[rng.Intn(len(keys))]
				} else {
					kb := make([]byte, 1+rng.Intn(40))
					rng.Read(kb)
					k = string(kb)
				}
				writes = append(writes, leafWrite{key: k, hash: v})
				merged[k] = v
			}
			checkApplied(t, fmt.Sprintf("iter %d (n=%d chunk=%d workers=%d)", iter, n, chunk, workers),
				mustApply(t, tr, changeSet(writes)), merged)
		})
	}
}

// Writes that leave every digest as it was — no inserts, same values — return
// the tree itself: nothing is copied and nothing is hashed.
func TestLeafTreeApplyNoOpKeepsTheTree(t *testing.T) {
	h := func(s string) [32]byte { return qcrypto.Hash32([]byte(s)) }
	tr := mustTree(t, map[string][32]byte{"b": h("b"), "d": h("d")})
	same := changeSet([]leafWrite{{key: "d", hash: h("d")}, {key: "b", hash: h("b")}})
	if got := mustApply(t, tr, same); got != tr {
		t.Fatal("a change set that changes nothing built a new tree")
	}
	if got := mustApply(t, tr, nil); got != tr {
		t.Fatal("an empty change set built a new tree")
	}
	moved := changeSet([]leafWrite{{key: "d", hash: h("d2")}})
	if got := mustApply(t, tr, moved); got == tr {
		t.Fatal("a real change returned the unchanged tree")
	}
}

func TestLeafTreeFind(t *testing.T) {
	h := func(s string) [32]byte { return qcrypto.Hash32([]byte(s)) }
	tr := mustTree(t, map[string][32]byte{"b": h("b"), "d": h("d"), "f": h("f")})
	for key, want := range map[string]struct {
		i  int
		ok bool
	}{"a": {0, false}, "b": {0, true}, "c": {1, false}, "d": {1, true}, "f": {2, true}, "g": {3, false}} {
		i, ok := tr.find(key)
		if i != want.i || ok != want.ok {
			t.Fatalf("find(%q) = (%d,%v), want (%d,%v)", key, i, ok, want.i, want.ok)
		}
	}
}

// A load that meets keys out of order must refuse: the root would silently
// cover a different ordering than every other node's.
func TestLeafTreeBuilderRefusesUnsortedKeys(t *testing.T) {
	var b leafTreeBuilder
	if err := b.add([]byte("b"), [32]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := b.add([]byte("a"), [32]byte{}); err == nil {
		t.Fatal("a key sorting before its predecessor was accepted")
	}
	var dup leafTreeBuilder
	if err := dup.add([]byte("k"), [32]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := dup.add([]byte("k"), [32]byte{}); err == nil {
		t.Fatal("a repeated key was accepted")
	}
}

// The chunk size nodes ship with, on trees of several chunks: every tail
// length around the chunk boundaries and more workers than some trees have
// chunks.
func TestLeafTreeRootMatchesReferenceAtShippingChunk(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for _, n := range []int{4095, 4096, 4097, 8191, 8192, 8193, 3*4096 + 1, 5*4096 - 1, 65535, 65536, 65537, 100003} {
		idx := randIndex(rng, n)
		want := merkleRoot(idx)
		for _, workers := range []int{1, 2, 3, 4, 7, 8} {
			withRootConfig(t, 4096, workers, func() {
				if got := mustTree(t, idx).root; got != want {
					t.Fatalf("n=%d workers=%d: %x, want %x", n, workers, got[:8], want[:8])
				}
			})
		}
	}
}

// Change sets applied to a tree of several shipping-size chunks — inserts
// first, last, adjacent and next to an update — and to the tree that results,
// as a promoted tree becomes the next block's base.
func TestLeafTreeApplyAtShippingChunk(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	base := randIndex(rng, 20000)
	keys := sortedKeys(base)
	withRootConfig(t, 4096, 4, func() {
		tr := mustTree(t, base)
		for iter := 0; iter < 25; iter++ {
			merged := make(map[string][32]byte, len(base)+64)
			for k, v := range base {
				merged[k] = v
			}
			var writes []leafWrite
			set := func(k string) {
				var v [32]byte
				rng.Read(v[:])
				writes = append(writes, leafWrite{key: k, hash: v})
				merged[k] = v
			}
			set("\x00")                 // sorts first
			set("\xff\xff\xff\xff\xff") // sorts last
			k := keys[rng.Intn(len(keys))]
			set(k)           // update
			set(k + "\x00")  // insert right after it
			set(k + "\x00a") // and another next to that
			for c := rng.Intn(40); c > 0; c-- {
				if rng.Intn(2) == 0 {
					set(keys[rng.Intn(len(keys))])
				} else {
					kb := make([]byte, 1+rng.Intn(40))
					rng.Read(kb)
					set(string(kb))
				}
			}
			got := mustApply(t, tr, changeSet(writes))
			checkApplied(t, fmt.Sprintf("iter %d", iter), got, merged)

			var next []leafWrite
			for c := 0; c < 5; c++ {
				kb := make([]byte, 1+rng.Intn(10))
				rng.Read(kb)
				var v [32]byte
				rng.Read(v[:])
				next = append(next, leafWrite{key: string(kb), hash: v})
				merged[string(kb)] = v
			}
			checkApplied(t, fmt.Sprintf("iter %d, next block", iter), mustApply(t, got, changeSet(next)), merged)
		}
	})
}

// Chunks only reduce exactly when they are powers of two; any other size must
// fall back to the sequential reduction rather than fork the root.
func TestLeafTreeRootIgnoresChunkSizesThatAreNotPowersOfTwo(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, n := range []int{5, 7, 13, 25, 37, 50, 97} {
		idx := randIndex(rng, n)
		want := merkleRoot(idx)
		for _, chunk := range []int{3, 6, 12} {
			withRootConfig(t, chunk, 4, func() {
				if got := mustTree(t, idx).root; got != want {
					t.Fatalf("n=%d chunk=%d: %x, want %x", n, chunk, got[:8], want[:8])
				}
			})
		}
	}
}
