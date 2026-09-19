package objects

import (
	"bytes"
	"sync"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

func newCachedSender(t *testing.T, priv *qcrypto.PrivateKey, seed byte, height uint64) *ObjectSender {
	t.Helper()
	send, err := NewObjectSender(bytes.Repeat([]byte{seed}, 64), ObjectDescriptor{
		Anchor:     Anchor{Kind: "block", ID: [32]byte{seed}, Height: height},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return send
}

func TestCachePutGetAndEviction(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	c := NewCache(2)
	if _, err := c.Get(make([]byte, 32)); err == nil {
		t.Fatal("empty cache served an object")
	}
	if _, err := c.Get([]byte{1}); err == nil {
		t.Fatal("short descriptor id accepted")
	}

	first := newCachedSender(t, priv, 1, 1)
	second := newCachedSender(t, priv, 2, 2)
	third := newCachedSender(t, priv, 3, 3)
	c.Put(first)
	c.Put(second)

	firstID := first.Manifest().DescriptorID()
	secondID := second.Manifest().DescriptorID()
	thirdID := third.Manifest().DescriptorID()

	got, err := c.Get(firstID[:])
	if err != nil {
		t.Fatalf("cached object lost: %v", err)
	}
	if got.Manifest().Anchor.Height != 1 {
		t.Fatal("wrong object returned")
	}

	c.Put(third) // evicts first (FIFO)
	if _, err := c.Get(firstID[:]); err == nil {
		t.Fatal("evicted object still served")
	}
	if _, err := c.Get(secondID[:]); err != nil {
		t.Fatalf("live object wrongly evicted: %v", err)
	}
	if _, err := c.Get(thirdID[:]); err != nil {
		t.Fatalf("newest object not served: %v", err)
	}

	// Re-Put with the same descriptor replaces in place without evicting:
	// identical anchor fields, so the descriptor ID is identical.
	replacement := newCachedSender(t, priv, 2, 2)
	c.Put(replacement)
	got, err = c.Get(secondID[:])
	if err != nil {
		t.Fatal(err)
	}
	if got != replacement {
		t.Fatal("same-descriptor Put did not replace")
	}
	if _, err := c.Get(thirdID[:]); err != nil {
		t.Fatalf("same-descriptor Put evicted a live entry: %v", err)
	}
	if len(c.order) != 2 {
		t.Fatalf("cache grew to %d entries on a replace, want 2", len(c.order))
	}
}

// The cache is shared by concurrent sealed handlers (batch Put vs symbol
// Get); run the race detector over a mixed load.
func TestCacheConcurrentPutGet(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	c := NewCache(256) // 200 distinct entries across the goroutines: no eviction
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed byte) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				send := newCachedSender(t, priv, seed+byte(i), uint64(i))
				c.Put(send)
				descID := send.Manifest().DescriptorID()
				if _, err := c.Get(descID[:]); err != nil {
					t.Errorf("concurrent get lost its own put: %v", err)
					return
				}
				c.Len()
			}
		}(byte(g))
	}
	wg.Wait()
}
