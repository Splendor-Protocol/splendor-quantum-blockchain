package objects

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
	"github.com/splendor-protocol/splendor-quantum-blockchain/network/coding"
)

func testObject(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}

func transferDesc(codec string, object []byte, symbolSize uint32) Descriptor {
	return Descriptor{
		Anchor:     Anchor{Kind: "block", ID: [32]byte{9, 9, 9}, Height: 7},
		Codec:      codec,
		ObjectSize: uint64(len(object)),
		SymbolSize: symbolSize,
	}
}

func mustSender(t *testing.T, object []byte, codec coding.Codec, repair int, priv *qcrypto.PrivateKey) *Sender {
	t.Helper()
	s, err := NewSender(object, transferDesc(codec.Name(), object, 64), codec, repair, priv, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func deliverAll(t *testing.T, recv *Receiver, send *Sender) {
	t.Helper()
	for i := 0; i < send.SymbolCount(); i++ {
		if recv.Ready() {
			return
		}
		p, err := send.Proof(i)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := recv.AbsorbProof(p); err != nil {
			t.Fatalf("absorb proof %d: %v", i, err)
		}
	}
}

func TestTransferRoundTripAllCodecs(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(31))
	// 1000 is not a multiple of 64, so the last symbol is padded.
	object := testObject(rng, 1000)
	for _, codec := range []coding.Codec{
		coding.NewUncodedCodec(),
		coding.NewErasureCodec(),
		coding.NewRLNCCodec(),
	} {
		t.Run(codec.Name(), func(t *testing.T) {
			send := mustSender(t, object, codec, 8, priv)
			recv, err := NewReceiver(send.Manifest(), DefaultLimits(), AllowSignerBytes(send.Manifest().SignerPubKey))
			if err != nil {
				t.Fatal(err)
			}
			deliverAll(t, recv, send)
			if !recv.Ready() {
				t.Fatalf("not ready after %d proofs, rank %d", send.SymbolCount(), recv.Rank())
			}
			got, err := recv.Decode()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, object) {
				t.Fatal("decoded object does not match source")
			}
		})
	}
}

func TestTransferRecoversFromSystematicLossWithRepair(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(32))
	object := testObject(rng, 16*64)
	codec := coding.NewRLNCCodec()
	send := mustSender(t, object, codec, 12, priv)
	recv, err := NewReceiver(send.Manifest(), DefaultLimits(), AllowSignerBytes(send.Manifest().SignerPubKey))
	if err != nil {
		t.Fatal(err)
	}
	src := int(send.Manifest().Descriptor.SourceCount)
	// Drop every third systematic leaf; fill rank from the finite repair set.
	for i := 0; i < send.SymbolCount(); i++ {
		if i < src && i%3 == 0 {
			continue
		}
		p, err := send.Proof(i)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := recv.AbsorbProof(p); err != nil {
			t.Fatal(err)
		}
		if recv.Ready() {
			break
		}
	}
	if !recv.Ready() {
		t.Fatalf("rank %d after loss+repair, want %d", recv.Rank(), src)
	}
	got, err := recv.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, object) {
		t.Fatal("loss+repair reconstructed the wrong object")
	}
}

func TestAbsorbProofRejectsTamperingBeforeDecode(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(33))
	object := testObject(rng, 8*64)
	send := mustSender(t, object, coding.NewRLNCCodec(), 4, priv)
	recv, err := NewReceiver(send.Manifest(), DefaultLimits(), AllowSignerBytes(send.Manifest().SignerPubKey))
	if err != nil {
		t.Fatal(err)
	}
	proof, err := send.Proof(0)
	if err != nil {
		t.Fatal(err)
	}

	tampered := proof
	tampered.Symbol.Payload = append([]byte(nil), proof.Symbol.Payload...)
	tampered.Symbol.Payload[0] ^= 0xff
	if _, err := recv.AbsorbProof(tampered); !errors.Is(err, ErrUnauthenticatedSymbol) {
		t.Fatalf("tampered payload = %v, want ErrUnauthenticatedSymbol", err)
	}
	if recv.Rank() != 0 {
		t.Fatalf("rank = %d after a failed proof: decoder was touched", recv.Rank())
	}

	badIdx := proof
	badIdx.Symbol.Index = int(send.Manifest().CodebookSize) + 1
	if _, err := recv.AbsorbProof(badIdx); !errors.Is(err, ErrUnauthenticatedSymbol) {
		t.Fatalf("out-of-range index = %v, want ErrUnauthenticatedSymbol", err)
	}
	if recv.Rank() != 0 {
		t.Fatal("out-of-range index increased rank")
	}
}

func TestAbsorbProofRejectsForeignCodebook(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(34))
	a := testObject(rng, 4*64)
	b := testObject(rng, 4*64)
	sendA := mustSender(t, a, coding.NewRLNCCodec(), 2, priv)
	sendB := mustSender(t, b, coding.NewRLNCCodec(), 2, priv)
	recv, err := NewReceiver(sendA.Manifest(), DefaultLimits(), AllowSignerBytes(sendA.Manifest().SignerPubKey))
	if err != nil {
		t.Fatal(err)
	}
	proofB, err := sendB.Proof(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recv.AbsorbProof(proofB); !errors.Is(err, ErrUnauthenticatedSymbol) {
		t.Fatalf("foreign proof = %v, want ErrUnauthenticatedSymbol", err)
	}
	if recv.Rank() != 0 {
		t.Fatal("a proof from another codebook increased rank")
	}
}

func TestNewReceiverRefusesUnauthorizedAndNilAuthorizer(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(35))
	object := testObject(rng, 128)
	send := mustSender(t, object, coding.NewUncodedCodec(), 0, priv)

	if _, err := NewReceiver(send.Manifest(), DefaultLimits(), nil); !errors.Is(err, ErrNilAuthorizer) {
		t.Fatalf("nil authorizer = %v, want ErrNilAuthorizer", err)
	}
	if _, err := NewReceiver(send.Manifest(), DefaultLimits(), AllowSignerBytes(otherPub.Bytes())); !errors.Is(err, ErrUnauthorizedSigner) {
		t.Fatalf("wrong signer = %v, want ErrUnauthorizedSigner", err)
	}
}

func TestDecodeBeforeReadyFails(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(36))
	object := testObject(rng, 4*64)
	send := mustSender(t, object, coding.NewRLNCCodec(), 2, priv)
	recv, err := NewReceiver(send.Manifest(), DefaultLimits(), AllowSignerBytes(send.Manifest().SignerPubKey))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recv.Decode(); !errors.Is(err, ErrReceiverNotReady) {
		t.Fatalf("empty decode = %v, want ErrReceiverNotReady", err)
	}
	p, err := send.Proof(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recv.AbsorbProof(p); err != nil {
		t.Fatal(err)
	}
	if _, err := recv.Decode(); !errors.Is(err, ErrReceiverNotReady) {
		t.Fatalf("partial decode = %v, want ErrReceiverNotReady", err)
	}
}

func TestNewSenderFillsAndChecksDescriptor(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	object := bytes.Repeat([]byte{1}, 100)
	codec := coding.NewUncodedCodec()

	// Codec name filled from the encoder when omitted.
	s, err := NewSender(object, Descriptor{
		Anchor:     Anchor{Kind: "snapshot", ID: [32]byte{1}, Height: 3},
		SymbolSize: 32,
	}, codec, 0, priv, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	m := s.Manifest()
	if m.Descriptor.Codec != codec.Name() {
		t.Fatalf("codec = %q, want %q", m.Descriptor.Codec, codec.Name())
	}
	if m.Descriptor.ObjectSize != 100 || m.Descriptor.SourceCount != 4 {
		t.Fatalf("filled sizes = %+v", m.Descriptor)
	}

	if _, err := NewSender(object, Descriptor{
		Anchor:     Anchor{Kind: "snapshot", ID: [32]byte{1}},
		Codec:      "rlnc-gf256-v1",
		SymbolSize: 32,
	}, codec, 0, priv, DefaultLimits()); err == nil {
		t.Fatal("mismatched codec name accepted")
	}
	if _, err := NewSender(object, Descriptor{
		Anchor:     Anchor{Kind: "snapshot", ID: [32]byte{1}},
		ObjectSize: 99,
		SymbolSize: 32,
	}, codec, 0, priv, DefaultLimits()); err == nil {
		t.Fatal("mismatched object size accepted")
	}
}

func TestSplitJoinPaddingRoundTrip(t *testing.T) {
	object := []byte("hello world") // 11 bytes
	syms, err := Split(object, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 2 || len(syms[0]) != 8 || len(syms[1]) != 8 {
		t.Fatalf("split shape = %d symbols", len(syms))
	}
	got, err := Join(syms, uint64(len(object)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, object) {
		t.Fatalf("join = %q, want %q", got, object)
	}
	syms[1][7] = 1 // corrupt padding
	if _, err := Join(syms, uint64(len(object))); err == nil {
		t.Fatal("nonzero padding accepted")
	}
}

func TestReceiverAcceptsWireRoundTrippedManifestAndProof(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(37))
	object := testObject(rng, 3*64)
	send := mustSender(t, object, coding.NewErasureCodec(), 2, priv)

	mFrame, err := EncodeManifest(send.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	m, err := DecodeManifest(mFrame)
	if err != nil {
		t.Fatal(err)
	}
	recv, err := NewReceiver(m, DefaultLimits(), AllowSignerBytes(m.SignerPubKey))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < send.SymbolCount(); i++ {
		p, err := send.Proof(i)
		if err != nil {
			t.Fatal(err)
		}
		pFrame, err := EncodeSymbolProof(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeSymbolProof(pFrame)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := recv.AbsorbProof(got); err != nil {
			t.Fatal(err)
		}
		if recv.Ready() {
			break
		}
	}
	decoded, err := recv.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, object) {
		t.Fatal("wire-framed transfer reconstructed the wrong object")
	}
}

func TestUncodedCannotRecoverDroppedSystematic(t *testing.T) {
	_, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(38))
	object := testObject(rng, 4*64)
	send := mustSender(t, object, coding.NewUncodedCodec(), 8, priv)
	if send.SymbolCount() != int(send.Manifest().Descriptor.SourceCount) {
		t.Fatalf("uncoded codebook size %d, want systematic-only %d",
			send.SymbolCount(), send.Manifest().Descriptor.SourceCount)
	}
	recv, err := NewReceiver(send.Manifest(), DefaultLimits(), AllowSignerBytes(send.Manifest().SignerPubKey))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < send.SymbolCount(); i++ { // drop index 0
		p, err := send.Proof(i)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := recv.AbsorbProof(p); err != nil {
			t.Fatal(err)
		}
	}
	if recv.Ready() {
		t.Fatal("uncoded transfer became ready after dropping a systematic symbol")
	}
}
