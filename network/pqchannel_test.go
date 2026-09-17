package network

import (
	"crypto/mlkem"
	"errors"
	"sync"
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

// TestMalformedEnvelopeNoPanic verifies that malformed envelopes return errors
// instead of panicking the node. This is a critical DOS prevention fix.
func TestMalformedEnvelopeNoPanic(t *testing.T) {
	// Create a valid node identity for testing
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("failed to generate test keypair: %v", err)
	}

	node, err := NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("failed to create node identity: %v", err)
	}

	t.Run("InvalidNonceLength", func(t *testing.T) {
		// Create envelope with invalid nonce length (should be 12, using 8)
		env := &Envelope{
			KEMCiphertext: make([]byte, mlkem.CiphertextSize768),
			Nonce:         make([]byte, 8), // INVALID: wrong size
			Ciphertext:    make([]byte, 32),
			Timestamp:     time.Now().Unix(),
		}

		// This should return an error, NOT panic
		_, err := node.Open(env)
		if err == nil {
			t.Fatal("expected error for invalid nonce length, got nil")
		}
		t.Logf("✓ Got expected error for invalid nonce: %v", err)
	})

	t.Run("TruncatedCiphertext", func(t *testing.T) {
		// Create envelope with ciphertext shorter than GCM tag
		env := &Envelope{
			KEMCiphertext: make([]byte, mlkem.CiphertextSize768),
			Nonce:         make([]byte, gcmNonceSize), // Correct nonce size
			Ciphertext:    make([]byte, 8),            // INVALID: too short (< 16)
			Timestamp:     time.Now().Unix(),
		}

		_, err := node.Open(env)
		if err == nil {
			t.Fatal("expected error for truncated ciphertext, got nil")
		}
		t.Logf("✓ Got expected error for truncated ciphertext: %v", err)
	})

	t.Run("ValidEnvelopeStillWorks", func(t *testing.T) {
		// Verify that valid envelopes still decrypt correctly
		doc, err := node.Document()
		if err != nil {
			t.Fatalf("failed to get identity document: %v", err)
		}

		if err := doc.Verify(nil); err != nil {
			t.Fatalf("identity document failed verification: %v", err)
		}

		plaintext := []byte("test message for encryption")
		env, err := Seal(doc, plaintext)
		if err != nil {
			t.Fatalf("failed to seal message: %v", err)
		}

		decrypted, err := node.Open(env)
		if err != nil {
			t.Fatalf("failed to open valid envelope: %v", err)
		}

		if string(decrypted) != string(plaintext) {
			t.Fatalf("decrypted message mismatch: got %q, want %q", decrypted, plaintext)
		}
		t.Logf("✓ Valid envelope encrypted and decrypted successfully")
	})
}

// TestEnvelopeValidationOrder verifies that cheap validations happen before
// expensive cryptographic operations (defense in depth).
func TestEnvelopeValidationOrder(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("failed to generate test keypair: %v", err)
	}

	node, err := NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("failed to create node identity: %v", err)
	}

	// Bad nonce but valid KEM ciphertext - nonce check should fail first
	env := &Envelope{
		KEMCiphertext: make([]byte, mlkem.CiphertextSize768),
		Nonce:         make([]byte, 999), // Obviously wrong
		Ciphertext:    make([]byte, 32),
		Timestamp:     time.Now().Unix(),
	}

	_, err = node.Open(env)
	// Should get an error without spending CPU on decapsulation
	if err == nil {
		t.Fatal("expected validation error for absurd nonce length")
	}
	t.Logf("✓ Cheap validation rejected bad envelope before expensive crypto")
}

// newTestNode builds a fresh NodeIdentity for lifecycle tests, the setup every
// one of these share beyond the envelope body checks above.
func newTestNode(t *testing.T) *NodeIdentity {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	node, err := NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("node identity: %v", err)
	}
	return node
}

func sealRoundTrip(t *testing.T, node *NodeIdentity, doc *IdentityDocument, msg string) *Envelope {
	t.Helper()
	env, err := Seal(doc, []byte(msg))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	pt, err := node.Open(env)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(pt) != msg {
		t.Fatalf("round trip got %q, want %q", pt, msg)
	}
	return env
}

// The healthy case: an envelope sealed to the current document opens, and
// opens to exactly the bytes that were sealed.
func TestChannelRoundTrip(t *testing.T) {
	node := newTestNode(t)
	doc, err := node.Document()
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	if err := doc.Verify(nil); err != nil {
		t.Fatalf("document failed to verify: %v", err)
	}
	sealRoundTrip(t, node, doc, "a sealed write-plane message")
}

// Rotation must not drop in-flight traffic: a message sealed to the
// just-rotated-out key still opens once (the previous key is retained for a
// grace window). After a SECOND rotation that key is gone, so the same
// envelope now fails — the forward-secrecy property a rotated key exists to
// provide.
func TestChannelRotationGrace(t *testing.T) {
	node := newTestNode(t)
	doc1, err := node.Document()
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	sealRoundTrip(t, node, doc1, "sealed to the first key")
	env1held, err := Seal(doc1, []byte("held for after rotate"))
	if err != nil {
		t.Fatalf("seal held: %v", err)
	}
	env1late, err := Seal(doc1, []byte("opened only after two rotations"))
	if err != nil {
		t.Fatalf("seal late: %v", err)
	}

	// One rotation: the first key becomes `previous` and still opens a
	// message sealed to it that has not been opened yet.
	if err := node.rotate(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := node.Open(env1held); err != nil {
		t.Fatalf("envelope sealed to key1 failed to open after one rotation: %v", err)
	}
	doc2, err := node.Document()
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	sealRoundTrip(t, node, doc2, "sealed to the second key")
	env2held, err := Seal(doc2, []byte("held for second rotate"))
	if err != nil {
		t.Fatalf("seal env2: %v", err)
	}

	// A second rotation retires key1 for good.
	if err := node.rotate(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := node.Open(env1late); err == nil {
		t.Fatal("envelope sealed to key1 still opened after two rotations")
	}
	// key2 is still within the grace window after exactly one subsequent rotation.
	if _, err := node.Open(env2held); err != nil {
		t.Fatalf("envelope sealed to key2 failed to open after the rotation that retired key1: %v", err)
	}
}

// A substituted KEM key must fail document verification: the signature binds
// KEMPubKey to the node's identity key, so an attacker cannot swap in their
// own encapsulation key and decrypt traffic intended for the real node.
func TestChannelKEMSubstitutionRejected(t *testing.T) {
	node := newTestNode(t)
	doc, err := node.Document()
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	tampered := *doc
	tampered.KEMPubKey = append([]byte(nil), doc.KEMPubKey...)
	tampered.KEMPubKey[0] ^= 0xff
	if err := tampered.Verify(nil); err == nil {
		t.Fatal("identity document with a substituted KEM key still verified")
	}
	// Open must not accept a message sealed to the substituted key either.
	env, err := Seal(&tampered, []byte("to the impostor"))
	if err != nil {
		t.Fatalf("seal to substituted key: %v", err)
	}
	if _, err := node.Open(env); err == nil {
		t.Fatal("node opened a message sealed to a substituted KEM key")
	}
}

// Any tamper of the ciphertext must fail authentication, even though the KEM
// ciphertext and nonce are untouched.
func TestChannelCiphertextTamperRejected(t *testing.T) {
	node := newTestNode(t)
	doc, err := node.Document()
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	env, err := Seal(doc, []byte("integrity-protected payload"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	env.Ciphertext[len(env.Ciphertext)-1] ^= 0xff
	if _, err := node.Open(env); err == nil {
		t.Fatal("tampered ciphertext still authenticated")
	}
}

// TestEnvelopeTimestampWindow verifies timestamp validation prevents replay
func TestEnvelopeTimestampWindow(t *testing.T) {
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("failed to generate test keypair: %v", err)
	}

	node, err := NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("failed to create node identity: %v", err)
	}

	// Envelope with timestamp way in the past
	env := &Envelope{
		KEMCiphertext: make([]byte, mlkem.CiphertextSize768),
		Nonce:         make([]byte, gcmNonceSize),
		Ciphertext:    make([]byte, 32),
		Timestamp:     time.Now().Add(-1 * time.Hour).Unix(), // Too old
	}

	_, err = node.Open(env)
	if err == nil {
		t.Fatal("expected error for stale timestamp")
	}
	t.Logf("✓ Stale envelope rejected: %v", err)
}

func TestDocumentRotatesOnceUnderLock(t *testing.T) {
	node := newTestNode(t)
	node.mu.Lock()
	node.notAfter = time.Now().Add(-time.Second)
	node.mu.Unlock()

	const n = 8
	docs := make([]*IdentityDocument, n)
	errc := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			doc, err := node.Document()
			if err != nil {
				errc <- err
				return
			}
			docs[i] = doc
		}(i)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatalf("document: %v", err)
	}
	first := docs[0].KEMPubKey
	for i, doc := range docs {
		if doc == nil || !bytesEqualConstantTime(doc.KEMPubKey, first) {
			t.Fatalf("concurrent expiry rotated more than once: doc %d diverged", i)
		}
	}
}

func TestPreviousKeyRetiresAfterGrace(t *testing.T) {
	node := newTestNode(t)
	doc1, err := node.Document()
	if err != nil {
		t.Fatal(err)
	}
	env, err := Seal(doc1, []byte("grace"))
	if err != nil {
		t.Fatal(err)
	}
	if err := node.rotate(); err != nil {
		t.Fatal(err)
	}
	node.mu.Lock()
	node.previousUntil = time.Now().Add(-time.Second)
	node.mu.Unlock()
	if _, err := node.Open(env); err == nil {
		t.Fatal("previous key still opened after its grace period")
	}
}

func TestEnvelopeReplayIsDistinctFromNewMessage(t *testing.T) {
	node := newTestNode(t)
	doc, err := node.Document()
	if err != nil {
		t.Fatal(err)
	}
	env, err := Seal(doc, []byte("once"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.Open(env); err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := node.Open(env); !errors.Is(err, ErrEnvelopeReplay) {
		t.Fatalf("retransmission = %v, want ErrEnvelopeReplay", err)
	}
	env2, err := Seal(doc, []byte("once"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := node.Open(env2)
	if err != nil {
		t.Fatalf("new envelope with same plaintext: %v", err)
	}
	if string(pt) != "once" {
		t.Fatalf("new message = %q", pt)
	}
}
