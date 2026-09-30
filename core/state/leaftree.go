package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

// rootChunkLeaves is the width of the aligned subtrees reduceRoot hashes in
// parallel. It must be a power of two of at least 2; anything else hashes
// sequentially. A variable so tests can drive small chunks.
var rootChunkLeaves = 4096

// rootWorkers is how many goroutines reduceRoot hashes chunks with.
var rootWorkers = func() int {
	return min(runtime.GOMAXPROCS(0), 8)
}

// leafTree is one immutable view of the committed leaf set: every leaf key in
// sorted order, each leaf's digest, and the Merkle root over them. Nothing
// mutates a tree once it is built — a commit installs a new one — so a reader
// holding a tree can walk it without the StateDB lock.
//
// Keys sit back to back in one arena indexed by offsets instead of in a slice
// of strings, so a tree holds no pointers for the garbage collector to trace;
// at a million leaves that is the difference between one object and a million.
type leafTree struct {
	arena []byte
	offs  []uint32   // key i is arena[offs[i]:offs[i+1]]; len(offs) == size()+1
	dig   [][32]byte // dig[i] is leafDigest(key i, value hash i)
	root  [32]byte
}

// size is the number of leaves. A nil tree (a simulation sandbox) has none.
func (t *leafTree) size() int {
	if t == nil {
		return 0
	}
	return len(t.dig)
}

// rootOrEmpty is the tree's root, or the empty-state root for a nil tree.
func (t *leafTree) rootOrEmpty() [32]byte {
	if t == nil {
		return stateEmptyRoot
	}
	return t.root
}

func (t *leafTree) keyBytes(i int) []byte { return t.arena[t.offs[i]:t.offs[i+1]] }

func (t *leafTree) key(i int) string { return string(t.keyBytes(i)) }

// find returns the index of key, or the index it would be inserted at and
// false when the tree does not hold it.
func (t *leafTree) find(key string) (int, bool) {
	n := t.size()
	i := sort.Search(n, func(i int) bool { return string(t.keyBytes(i)) >= key })
	return i, i < n && string(t.keyBytes(i)) == key
}

// leafTreeBuilder assembles a tree from leaves supplied in strictly increasing
// key order, which is the order LevelDB iterates in.
type leafTreeBuilder struct {
	arena []byte
	offs  []uint32
	dig   [][32]byte
}

func (b *leafTreeBuilder) add(key []byte, d [32]byte) error {
	if len(b.offs) == 0 {
		b.offs = append(b.offs, 0)
	}
	if n := len(b.dig); n > 0 {
		if prev := b.arena[b.offs[n-1]:]; bytes.Compare(prev, key) >= 0 {
			return fmt.Errorf("state: leaf key %q does not sort after %q", key, prev)
		}
	}
	// Offsets are uint32, so all keys together are capped at 4 GiB — some 60
	// million account leaves. Every node reaches the cap at the same block and
	// stops there, which is a halt rather than a fork; widen the offsets long
	// before the state gets near it.
	if uint64(len(b.arena))+uint64(len(key)) > math.MaxUint32 {
		return errors.New("state: leaf keys exceed the 4 GiB index arena")
	}
	b.arena = append(b.arena, key...)
	b.offs = append(b.offs, uint32(len(b.arena)))
	b.dig = append(b.dig, d)
	return nil
}

func (b *leafTreeBuilder) build() *leafTree {
	if len(b.offs) == 0 {
		b.offs = []uint32{0}
	}
	return &leafTree{arena: b.arena, offs: b.offs, dig: b.dig, root: reduceRoot(b.dig)}
}

// leafTreeFromIndex builds the tree over a leaf index (key -> value hash), as
// a snapshot import holds it.
func leafTreeFromIndex(index map[string][32]byte) (*leafTree, error) {
	keys := make([]string, 0, len(index))
	size := 0
	for k := range index {
		keys = append(keys, k)
		size += len(k)
	}
	sort.Strings(keys)
	b := leafTreeBuilder{
		arena: make([]byte, 0, size),
		offs:  make([]uint32, 0, len(keys)+1),
		dig:   make([][32]byte, 0, len(keys)),
	}
	for _, k := range keys {
		if err := b.add([]byte(k), leafDigest(k, index[k])); err != nil {
			return nil, err
		}
	}
	return b.build(), nil
}

// leafWrite is one value the pending layer would commit, in commit order.
type leafWrite struct {
	key  string
	data []byte
	hash [32]byte // SHA3-256 of data
}

// leafChange is one leaf a commit sets: its key and its new digest.
type leafChange struct {
	key string
	dig [32]byte
}

// changeSet reduces ordered writes to the change set a tree applies: one entry
// per key, sorted, the last write to a key winning as it does in the commit
// batch.
func changeSet(writes []leafWrite) []leafChange {
	if len(writes) == 0 {
		return nil
	}
	last := make(map[string]int, len(writes))
	for i, w := range writes {
		last[w.key] = i
	}
	out := make([]leafChange, 0, len(last))
	for k, i := range last {
		out = append(out, leafChange{key: k, dig: leafDigest(k, writes[i].hash)})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].key < out[b].key })
	return out
}

func equalChanges(a, b []leafChange) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// apply returns the tree with delta applied — each change replaces an
// existing leaf's digest or inserts a new leaf — and leaves t as it was. delta
// must be sorted with no repeated key, as changeSet returns it.
//
// The work is one pass over the leaf arrays plus reduceRoot. Leaves are
// addressed by position in key order, so a key inserted anywhere but the end
// shifts every pair after it; that is why the internal levels are rehashed in
// full rather than patched.
func (t *leafTree) apply(delta []leafChange) (*leafTree, error) {
	if len(delta) == 0 {
		return t, nil
	}
	n := t.size()
	pos := make([]int, len(delta))
	found := make([]bool, len(delta))
	inserts, insertBytes := 0, 0
	for i, c := range delta {
		pos[i], found[i] = t.find(c.key)
		if !found[i] {
			inserts++
			insertBytes += len(c.key)
		}
	}

	if inserts == 0 {
		unchanged := true
		for i, c := range delta {
			if t.dig[pos[i]] != c.dig {
				unchanged = false
				break
			}
		}
		if unchanged {
			return t, nil
		}
		// Same keys: share the key arena, replace digests in a copy.
		dig := make([][32]byte, n)
		copy(dig, t.dig)
		for i, c := range delta {
			dig[pos[i]] = c.dig
		}
		return &leafTree{arena: t.arena, offs: t.offs, dig: dig, root: reduceRoot(dig)}, nil
	}

	baseArena := 0
	if t != nil {
		baseArena = len(t.arena)
	}
	if uint64(baseArena)+uint64(insertBytes) > math.MaxUint32 { // see leafTreeBuilder.add
		return nil, errors.New("state: leaf keys exceed the 4 GiB index arena")
	}
	m := n + inserts
	arena := make([]byte, 0, baseArena+insertBytes)
	offs := make([]uint32, 1, m+1)
	dig := make([][32]byte, 0, m)

	// copyBase appends base leaves [a, b) unchanged.
	copyBase := func(a, b int) {
		if a >= b {
			return
		}
		start := uint32(len(arena))
		arena = append(arena, t.arena[t.offs[a]:t.offs[b]]...)
		for i := a; i < b; i++ {
			offs = append(offs, t.offs[i+1]-t.offs[a]+start)
		}
		dig = append(dig, t.dig[a:b]...)
	}
	addLeaf := func(key []byte, d [32]byte) {
		arena = append(arena, key...)
		offs = append(offs, uint32(len(arena)))
		dig = append(dig, d)
	}

	next := 0 // the first base leaf not yet copied
	for i, c := range delta {
		copyBase(next, pos[i])
		if found[i] {
			addLeaf(t.keyBytes(pos[i]), c.dig)
			next = pos[i] + 1
		} else {
			addLeaf([]byte(c.key), c.dig)
			next = pos[i]
		}
	}
	copyBase(next, n)
	return &leafTree{arena: arena, offs: offs, dig: dig, root: reduceRoot(dig)}, nil
}

// proofSteps walks leaf i up to the root, recording each sibling and the side
// it sits on. A carried odd node has no sibling and adds no step.
func (t *leafTree) proofSteps(ctx context.Context, i int) ([]MerkleStep, error) {
	level := t.dig
	var steps []MerkleStep
	idx := i
	for len(level) > 1 {
		next := make([][32]byte, 0, (len(level)+1)/2)
		for j := 0; j < len(level); j += 2 {
			if j&8191 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if j+1 == len(level) {
				next = append(next, level[j])
				break
			}
			left, right := level[j], level[j+1]
			next = append(next, internalHash(left, right))
			if idx == j {
				steps = append(steps, MerkleStep{Sibling: right, Left: false})
			} else if idx == j+1 {
				steps = append(steps, MerkleStep{Sibling: left, Left: true})
			}
		}
		idx /= 2
		level = next
	}
	return steps, nil
}

// reduceRoot is the Merkle root over leaf digests already in key order: pairs
// hash left to right and an odd last node is carried up unchanged, exactly as
// merkleRoot builds it.
//
// Large trees are hashed in parallel in aligned chunks of rootChunkLeaves
// leaves, and that is exact rather than an approximation. A full chunk starts
// at an even index on every level below its own root, so no pair straddles it
// and it never carries. The tail chunk is the last run on every level, so its
// carries are the tree's own. The level the chunks reduce to is therefore just
// their roots in order, and the rest is the ordinary reduction over those.
func reduceRoot(dig [][32]byte) [32]byte {
	n := len(dig)
	if n == 0 {
		return stateEmptyRoot
	}
	chunk, workers := rootChunkLeaves, rootWorkers()
	if workers < 2 || chunk < 2 || chunk&(chunk-1) != 0 || n <= chunk {
		return reduceInto(dig, make([][32]byte, (n+1)/2))
	}
	chunks := (n + chunk - 1) / chunk
	workers = min(workers, chunks)
	roots := make([][32]byte, chunks)
	var claim atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scratch := make([][32]byte, (chunk+1)/2)
			for {
				c := int(claim.Add(1)) - 1
				if c >= chunks {
					return
				}
				lo := c * chunk
				roots[c] = reduceInto(dig[lo:min(lo+chunk, n)], scratch)
			}
		}()
	}
	wg.Wait()
	return reduceInto(roots, make([][32]byte, (chunks+1)/2))
}

// reduceInto hashes level up to its root without modifying it, using scratch
// (at least (len(level)+1)/2 entries) as the working level.
func reduceInto(level, scratch [][32]byte) [32]byte {
	if len(level) == 1 {
		return level[0]
	}
	m := 0
	for i := 0; i+1 < len(level); i += 2 {
		scratch[m] = internalHash(level[i], level[i+1])
		m++
	}
	if len(level)%2 == 1 {
		scratch[m] = level[len(level)-1]
		m++
	}
	for m > 1 {
		j := 0
		for i := 0; i+1 < m; i += 2 {
			scratch[j] = internalHash(scratch[i], scratch[i+1])
			j++
		}
		if m%2 == 1 {
			scratch[j] = scratch[m-1]
			j++
		}
		m = j
	}
	return scratch[0]
}
