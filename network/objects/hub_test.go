package objects

import (
	"bytes"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

func TestHubServesCurrentAndRejectsUnknown(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	object := bytes.Repeat([]byte{3}, 128)
	send, err := NewObjectSender(object, ObjectDescriptor{
		Anchor:     Anchor{Kind: "block", ID: [32]byte{1}, Height: 1},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub()
	if _, err := h.ServeManifest(nil); err == nil {
		t.Fatal("empty hub served a manifest")
	}
	h.Put(send)
	frame, err := h.ServeManifest(nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := DecodeObjectManifest(frame)
	if err != nil {
		t.Fatal(err)
	}
	id := m.DescriptorID()
	again, err := h.ServeManifest(id[:])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame, again) {
		t.Fatal("current vs descriptor-id lookup returned different frames")
	}
	proof, err := h.ServeSymbol(id[:], 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSymbolProof(proof); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ServeSymbol(id[:], 0, uint32(send.SymbolCount(0))); err == nil {
		t.Fatal("out-of-range symbol served")
	}
	if _, err := h.ServeSymbol(id[:], 1, 0); err == nil {
		t.Fatal("out-of-range generation served")
	}
	var other [32]byte
	other[0] = 0xff
	if _, err := h.ServeManifest(other[:]); err == nil {
		t.Fatal("unknown descriptor id served")
	}
}

func TestHubPutReplacesPrevious(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewObjectSender(bytes.Repeat([]byte{1}, 64), ObjectDescriptor{
		Anchor:     Anchor{Kind: "block", ID: [32]byte{1}, Height: 1},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewObjectSender(bytes.Repeat([]byte{2}, 64), ObjectDescriptor{
		Anchor:     Anchor{Kind: "block", ID: [32]byte{2}, Height: 2},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub()
	h.Put(first)
	old := first.Manifest().DescriptorID()
	h.Put(second)

	// The current object is the new one: a joiner that names no id is always
	// offered the freshest state, which is the point of refreshing at all.
	cur := h.Current()
	if cur.Manifest().Anchor.Height != 2 {
		t.Fatal("current object was not replaced")
	}

	// The replaced object is NOT gone the instant it is replaced -- it stays
	// addressable by its own descriptor id so a fetch already under way can
	// finish. This assertion used to read the other way, and that is exactly
	// why a refresh tore in-flight transfers; see
	// TestHubDoesNotTearAnInFlightFetch.
	if _, err := h.ServeManifest(old[:]); err != nil {
		t.Fatalf("the replaced object became unreachable immediately: %v", err)
	}

	// It does not stay for ever. Past the retirement TTL it is unmapped and
	// its codebook can be collected.
	h.now = func() time.Time { return time.Now().Add(RetiredTTL + time.Second) }
	if _, err := h.ServeManifest(old[:]); err == nil {
		t.Fatal("a retired object was still served after its TTL expired")
	}
	if h.Current() == nil {
		t.Fatal("expiry swept the current object")
	}
}

// A refresh must not tear a transfer already in flight. A fetch is a manifest
// read followed by one sealed request per symbol, all addressed by the
// descriptor id read out of that manifest -- so deleting the previous object
// on Put failed every remaining symbol with "no coded object published",
// discarding a verified manifest and every symbol absorbed under it, and
// saying something untrue while doing it.
func TestHubDoesNotTearAnInFlightFetch(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	object := func(fill byte, height uint64) *ObjectSender {
		t.Helper()
		o, oerr := NewObjectSender(bytes.Repeat([]byte{fill}, 256), ObjectDescriptor{
			Anchor:     Anchor{Kind: "snapshot", ID: [32]byte{fill}, Height: height},
			SymbolSize: 64,
		}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
		if oerr != nil {
			t.Fatal(oerr)
		}
		return o
	}

	h := NewHub()
	first := object(1, 100)
	h.Put(first)

	// The fetcher reads the manifest and takes the first symbol.
	frame, err := h.ServeManifest(nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := DecodeObjectManifest(frame)
	if err != nil {
		t.Fatal(err)
	}
	id := m.DescriptorID()
	if _, err := h.ServeSymbol(id[:], 0, 0); err != nil {
		t.Fatalf("symbol 0 of the object the fetcher was handed: %v", err)
	}

	// The publisher refreshes mid-fetch.
	h.Put(object(2, 356))

	// The rest of the fetch must still complete against the manifest the
	// fetcher verified.
	for i := uint32(1); i < uint32(first.SymbolCount(0)); i++ {
		if _, err := h.ServeSymbol(id[:], 0, i); err != nil {
			t.Fatalf("symbol %d became unfetchable the moment a newer object was published: %v", i, err)
		}
	}
	if _, err := h.ServeManifest(id[:]); err != nil {
		t.Fatalf("the in-flight object's own manifest became unfetchable: %v", err)
	}
}

// Memory is bounded by the number of retired generations, not by how often a
// publisher refreshes: the oldest falls out as newer ones arrive.
func TestHubRetirementIsBounded(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub()
	ids := make([][32]byte, 0, 6)
	for i := byte(1); i <= 6; i++ {
		o, oerr := NewObjectSender(bytes.Repeat([]byte{i}, 64), ObjectDescriptor{
			Anchor:     Anchor{Kind: "block", ID: [32]byte{i}, Height: uint64(i)},
			SymbolSize: 64,
		}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
		if oerr != nil {
			t.Fatal(oerr)
		}
		ids = append(ids, o.Manifest().DescriptorID())
		h.Put(o)
	}
	if got := len(h.byID); got != maxRetiredObjects+1 {
		t.Fatalf("hub holds %d objects, want %d (current + %d retired)", got, maxRetiredObjects+1, maxRetiredObjects)
	}
	for i, id := range ids {
		_, err := h.ServeManifest(id[:])
		wantReachable := i >= len(ids)-(maxRetiredObjects+1)
		if wantReachable && err != nil {
			t.Fatalf("object %d should still be reachable: %v", i+1, err)
		}
		if !wantReachable && err == nil {
			t.Fatalf("object %d should have been evicted", i+1)
		}
	}
}

func TestHubClearDropsCurrent(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	send, err := NewObjectSender(bytes.Repeat([]byte{5}, 64), ObjectDescriptor{
		Anchor:     Anchor{Kind: "block", ID: [32]byte{3}, Height: 3},
		SymbolSize: 64,
	}, coding.NewUncodedCodec(), 0, priv, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub()
	h.Put(send)
	h.Clear()
	if h.Current() != nil {
		t.Fatal("cleared hub still holds a current object")
	}
	if _, err := h.ServeManifest(nil); err == nil {
		t.Fatal("cleared hub served a manifest")
	}
}
