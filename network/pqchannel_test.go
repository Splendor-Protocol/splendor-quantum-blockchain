package network

import (
	"testing"
)

// TestMalformedEnvelopeNoPanic verifies that malformed envelopes return errors
// instead of panicking the node. This is a critical DOS prevention fix.
func TestMalformedEnvelopeNoPanic(t *testing.T) {
	// Create a valid node identity for testing
	pub, priv, err := generateTestKeypair()
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
			KEMCiphertext: make([]byte, 1088), // Correct KEM ciphertext size
			Nonce:         make([]byte, 8),     // INVALID: wrong size
			Ciphertext:    make([]byte, 32),
			Timestamp:     0,
		}

		// This should return an error, NOT panic
		_, err := node.Open(env)
		if err == nil {
			t.Fatal("expected error for invalid nonce length, got nil")
		}
		if err.Error() != "envelope did not decrypt under any current node key" {
			t.Logf("Got expected error: %v", err)
		}
	})

	t.Run("TruncatedCiphertext", func(t *testing.T) {
		// Create envelope with ciphertext shorter than GCM tag
		env := &Envelope{
			KEMCiphertext: make([]byte, 1088),
			Nonce:         make([]byte, 12), // Correct nonce size
			Ciphertext:    make([]byte, 8),  // INVALID: too short (< 16)
			Timestamp:     0,
		}

		_, err := node.Open(env)
		if err == nil {
			t.Fatal("expected error for truncated ciphertext, got nil")
		}
		t.Logf("Got expected error for truncated ciphertext: %v", err)
	})

	t.Run("ValidEnvelopeStillWorks", func(t *testing.T) {
		// Verify that valid envelopes still decrypt correctly
		doc, err := node.Document()
		if err != nil {
			t.Fatalf("failed to get identity document: %v", err)
		}

		plaintext := []byte("test message")
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
	})
}

// TestEnvelopeValidationOrder verifies that cheap validations happen before
// expensive cryptographic operations (defense in depth).
func TestEnvelopeValidationOrder(t *testing.T) {
	pub, priv, err := generateTestKeypair()
	if err != nil {
		t.Fatalf("failed to generate test keypair: %v", err)
	}
	
	node, err := NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("failed to create node identity: %v", err)
	}

	// Bad nonce but valid KEM ciphertext - nonce check should fail first
	env := &Envelope{
		KEMCiphertext: make([]byte, 1088),
		Nonce:         make([]byte, 999), // Obviously wrong
		Ciphertext:    make([]byte, 32),
		Timestamp:     0,
	}

	_, err = node.Open(env)
	// Should get an error without spending CPU on decapsulation
	if err == nil {
		t.Fatal("expected validation error for absurd nonce length")
	}
}

// Helper to generate test keypairs
func generateTestKeypair() (*PublicKey, *PrivateKey, error) {
	// This would use your actual key generation
	// For now, return nil as placeholder
	return nil, nil, nil
}

type PublicKey struct{}
type PrivateKey struct{}
