package network

import (
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/objects"
)

// makeReceiver builds a real, verified coded-object receiver with a distinct
// descriptor ID for each anchor, so cache-key collisions and eviction can be
// exercised with honest values. One keypair is reused across anchors: the
// descriptor ID derives from the anchor, not the signer.
func makeReceiver(t *testing.T, pub *qcrypto.PublicKey, priv *qcrypto.PrivateKey, anchor [32]byte) (*objects.ObjectReceiver, [32]byte) {
	t.Helper()
	desc := objects.ObjectDescriptor{
		Anchor:     objects.Anchor{Kind: "snapshot", ID: anchor, Height: 1},
		Codec:      coding.NewUncodedCodec().Name(),
		SymbolSize: 64,
	}
	send, err := objects.NewObjectSender([]byte("test coded object payload"), desc, coding.NewUncodedCodec(), 1, priv, objects.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	recv, err := objects.NewObjectReceiver(send.Manifest(), objects.DefaultLimits(), objects.AllowObjectSignerBytes(pub.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	return recv, send.Manifest().DescriptorID()
}

func TestObjectProgressKeepsAndResumes(t *testing.T) {
	p := newObjectProgress()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	recv, descID := makeReceiver(t, pub, priv, [32]byte{1})
	p.keep("peer-a", descID, recv, 64)

	got := p.take("peer-a", descID)
	if got != recv {
		t.Fatal("take did not return the retained receiver")
	}
	if p.Resumes() != 1 {
		t.Fatalf("Resumes = %d, want 1", p.Resumes())
	}
	if p.take("peer-a", descID) != nil {
		t.Fatal("a second take for the same key must be empty: the entry was consumed")
	}
}

func TestObjectProgressKeysByPeerAndDescriptor(t *testing.T) {
	p := newObjectProgress()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	recvA, idA := makeReceiver(t, pub, priv, [32]byte{1})
	recvB, idB := makeReceiver(t, pub, priv, [32]byte{2})

	p.keep("peer-a", idA, recvA, 64)
	p.keep("peer-a", idB, recvB, 64)
	// Same descriptor, different peer: a distinct entry.
	p.keep("peer-b", idA, recvB, 64)

	if got := p.take("peer-a", idB); got != recvB {
		t.Fatal("peer-a/idB take did not return recvB")
	}
	if got := p.take("peer-b", idA); got != recvB {
		t.Fatal("peer-b/idA take did not return its entry")
	}
	if got := p.take("peer-a", idA); got != recvA {
		t.Fatal("peer-a/idA take did not return recvA")
	}
}

func TestObjectProgressEvictsOldest(t *testing.T) {
	p := newObjectProgress()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	n := maxObjectProgressReceivers + 1
	receivers := make([]*objects.ObjectReceiver, n)
	ids := make([][32]byte, n)
	for i := 0; i < n; i++ {
		receivers[i], ids[i] = makeReceiver(t, pub, priv, [32]byte{byte(i + 1)})
		p.keep("peer-a", ids[i], receivers[i], 64)
	}
	// The oldest entry (i=0) was evicted; the rest are retained.
	if p.take("peer-a", ids[0]) != nil {
		t.Fatal("the oldest entry should have been evicted")
	}
	for i := 1; i < n; i++ {
		if got := p.take("peer-a", ids[i]); got != receivers[i] {
			t.Fatalf("entry %d was not retained", i)
		}
	}
}

func TestObjectProgressRejectsOversized(t *testing.T) {
	p := newObjectProgress()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	recv, id := makeReceiver(t, pub, priv, [32]byte{9})
	p.keep("peer-a", id, recv, maxObjectProgressBytes+1)
	if p.take("peer-a", id) != nil {
		t.Fatal("an object larger than the whole budget must not be retained")
	}
}
