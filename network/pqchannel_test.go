package network

import (
	"bytes"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

func newTestIdentity(t *testing.T) *NodeIdentity {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	id, err := NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	return id
}

// A sealed message must round-trip, and must be opaque on the wire.
func TestSealOpenRoundTrip(t *testing.T) {
	id := newTestIdentity(t)
	doc, err := id.Document()
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	if err := doc.Verify(nil); err != nil {
		t.Fatalf("self-issued document failed verification: %v", err)
	}

	plaintext := []byte(`{"header":{"height":42},"secret":"pre-broadcast tx"}`)
	env, err := Seal(doc, plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// The payload must not appear anywhere in the transmitted envelope.
	wire, err := MarshalEnvelope(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(wire, []byte("pre-broadcast tx")) {
		t.Fatal("plaintext leaked into the sealed envelope")
	}

	got, err := id.Open(env)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: got %q", got)
	}
}

// An envelope sealed to one node must not open under another node's key.
func TestSealedEnvelopeIsNodeSpecific(t *testing.T) {
	alice := newTestIdentity(t)
	bob := newTestIdentity(t)

	doc, _ := alice.Document()
	env, err := Seal(doc, []byte("for alice only"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := bob.Open(env); err == nil {
		t.Fatal("envelope sealed to alice opened under bob's key")
	}
}

// A machine-in-the-middle swapping in its own ML-KEM key must be detected: the
// identity signature is what binds the KEM key to the node identity.
func TestIdentityDocumentRejectsSubstitutedKEMKey(t *testing.T) {
	honest := newTestIdentity(t)
	attacker := newTestIdentity(t)

	doc, _ := honest.Document()
	attackerDoc, _ := attacker.Document()

	// Attacker keeps the honest node's advertised identity but swaps the KEM key
	// for one it can decapsulate.
	forged := *doc
	forged.KEMPubKey = attackerDoc.KEMPubKey

	if err := forged.Verify(nil); err == nil {
		t.Fatal("substituted KEM key passed verification — channel is MITM-able")
	}
}

// Pinning must reject a document signed by a different, otherwise-valid node.
func TestIdentityPinning(t *testing.T) {
	real := newTestIdentity(t)
	impostor := newTestIdentity(t)

	realDoc, _ := real.Document()
	impostorDoc, _ := impostor.Document()

	if err := impostorDoc.Verify(realDoc.NodePubKey); err == nil {
		t.Fatal("impostor document accepted against a pinned key")
	}
	if err := realDoc.Verify(realDoc.NodePubKey); err != nil {
		t.Fatalf("genuine document rejected against its own pinned key: %v", err)
	}
}

// Tampering with any authenticated field must break the AEAD tag.
func TestEnvelopeTamperDetection(t *testing.T) {
	id := newTestIdentity(t)
	doc, _ := id.Document()
	env, err := Seal(doc, []byte("block body"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	t.Run("ciphertext", func(t *testing.T) {
		tampered := *env
		tampered.Ciphertext = append([]byte(nil), env.Ciphertext...)
		tampered.Ciphertext[0] ^= 0xFF
		if _, err := id.Open(&tampered); err == nil {
			t.Fatal("modified ciphertext opened successfully")
		}
	})

	t.Run("timestamp", func(t *testing.T) {
		tampered := *env
		tampered.Timestamp = env.Timestamp - 1
		if _, err := id.Open(&tampered); err == nil {
			t.Fatal("modified timestamp opened successfully")
		}
	})
}

// A captured envelope must not replay outside the freshness window.
func TestEnvelopeReplayWindow(t *testing.T) {
	id := newTestIdentity(t)
	doc, _ := id.Document()
	env, err := Seal(doc, []byte("replay me"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	env.Timestamp = time.Now().Add(-2 * maxEnvelopeAge).Unix()
	if _, err := id.Open(env); err == nil {
		t.Fatal("stale envelope was accepted")
	}
}

// After rotation the previous key must still open in-flight messages, but a
// key from two rotations ago must not.
func TestKEMKeyRotation(t *testing.T) {
	id := newTestIdentity(t)
	doc, _ := id.Document()
	env, err := Seal(doc, []byte("in flight"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if err := id.rotate(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := id.Open(env); err != nil {
		t.Fatalf("in-flight message dropped by a single rotation: %v", err)
	}

	if err := id.rotate(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := id.Open(env); err == nil {
		t.Fatal("message still opened two rotations later — old keys are not being retired")
	}
}
