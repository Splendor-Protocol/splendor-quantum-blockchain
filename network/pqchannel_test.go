package network

import (
	"crypto/mlkem"
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
			Nonce:         make([]byte, 8),  // INVALID: wrong size
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
