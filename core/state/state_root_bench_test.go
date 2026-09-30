package state

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// benchLeafIndex returns n account-shaped leaves ("acc:SQ:" + 64 hex, the
// shape of the testnet heartbeat's fresh recipients) with random value hashes.
func benchLeafIndex(rng *rand.Rand, n int) map[string][32]byte {
	idx := make(map[string][32]byte, n)
	var a [32]byte
	for len(idx) < n {
		rng.Read(a[:])
		var v [32]byte
		rng.Read(v[:])
		idx[prefixAccount+"SQ:"+hex.EncodeToString(a[:])] = v
	}
	return idx
}

// BenchmarkStateRoot compares one root the old way (merkleRoot: sort every
// key, hash every leaf, then every level) with the tree the state DB now keeps,
// at the testnet's size and at a million leaves. Run on a Mac with -tags purego
// to hash with the same generic Keccak a Linux arm64 node uses.
//
//	reference   the old per-root cost; the old node paid it twice a block
//	heartbeat   a block like testnet's: two balances change, one fresh address
//	insertAt0   the worst single insert: the new key sorts first
//	updateOnly  two balances change, nobody new
//	proof       one account proof from the cached digests
func BenchmarkStateRoot(b *testing.B) {
	if testing.Short() {
		b.Skip("builds trees of up to a million leaves")
	}
	for _, n := range []int{370_000, 1_000_000} {
		rng := rand.New(rand.NewSource(int64(n)))
		idx := benchLeafIndex(rng, n)
		tree, err := leafTreeFromIndex(idx)
		if err != nil {
			b.Fatal(err)
		}
		change := func(key string) leafChange {
			var v [32]byte
			rng.Read(v[:])
			return leafChange{key: key, dig: leafDigest(key, v)}
		}
		existing := func() string { return tree.key(rng.Intn(n)) }
		fresh := func() string {
			var a [32]byte
			rng.Read(a[:])
			return prefixAccount + "SQ:" + hex.EncodeToString(a[:])
		}
		sorted := func(cs ...leafChange) []leafChange {
			sort.Slice(cs, func(i, j int) bool { return cs[i].key < cs[j].key })
			out := cs[:0]
			for _, c := range cs {
				if len(out) > 0 && out[len(out)-1].key == c.key {
					continue
				}
				out = append(out, c)
			}
			return out
		}
		apply := func(b *testing.B, delta func() []leafChange) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				d := delta()
				b.StartTimer()
				if _, err := tree.apply(d); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
			}
		}

		b.Run(fmt.Sprintf("leaves=%d/reference", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				merkleRoot(idx)
			}
		})
		b.Run(fmt.Sprintf("leaves=%d/heartbeat", n), func(b *testing.B) {
			b.StopTimer()
			apply(b, func() []leafChange { return sorted(change(existing()), change(existing()), change(fresh())) })
		})
		b.Run(fmt.Sprintf("leaves=%d/insertAt0", n), func(b *testing.B) {
			b.StopTimer()
			apply(b, func() []leafChange { return sorted(change("\x00")) })
		})
		b.Run(fmt.Sprintf("leaves=%d/updateOnly", n), func(b *testing.B) {
			b.StopTimer()
			apply(b, func() []leafChange { return sorted(change(existing()), change(existing())) })
		})
		b.Run(fmt.Sprintf("leaves=%d/proof", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := tree.proofSteps(context.Background(), n/2); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
