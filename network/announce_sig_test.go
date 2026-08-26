package network

import (
	"testing"
	"time"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

func testIdentity(t *testing.T) *NodeIdentity {
	t.Helper()
	pub, priv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	id, err := NewNodeIdentity(pub, priv)
	if err != nil {
		t.Fatalf("new node identity: %v", err)
	}
	return id
}

func TestSignedAnnounceRoundTrip(t *testing.T) {
	id := testIdentity(t)
	self := PeerInfo{Addr: "https://node-a.example:8080", Height: 42, Version: "v0.4.3"}

	sa, err := SignAnnounce(id, self)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := sa.Verify(NewAnnounceNonceCache(), time.Now()); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestSignedAnnounceRejectsForeignKey(t *testing.T) {
	id := testIdentity(t)
	other := testIdentity(t)
	otherPub, _ := other.SigningKeys()

	sa, err := SignAnnounce(id, PeerInfo{Addr: "https://node-a.example:8080"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Present someone else's key for a signature they did not make. This is the
	// whole point of the change: a signature names one node, where the shared
	// token named none.
	sa.NodePubKey = otherPub.Bytes()

	if err := sa.Verify(NewAnnounceNonceCache(), time.Now()); err == nil {
		t.Fatal("expected verification to fail with a substituted public key")
	}
}

func TestSignedAnnounceRejectsTamperedAddr(t *testing.T) {
	id := testIdentity(t)
	sa, err := SignAnnounce(id, PeerInfo{Addr: "https://node-a.example:8080", Height: 7})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sa.Peer.Addr = "https://attacker.example:8080"

	if err := sa.Verify(NewAnnounceNonceCache(), time.Now()); err == nil {
		t.Fatal("expected verification to fail after the announced address was changed")
	}
}

func TestSignedAnnounceRejectsReplay(t *testing.T) {
	id := testIdentity(t)
	cache := NewAnnounceNonceCache()
	sa, err := SignAnnounce(id, PeerInfo{Addr: "https://node-a.example:8080"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if err := sa.Verify(cache, time.Now()); err != nil {
		t.Fatalf("first delivery should verify: %v", err)
	}
	if err := sa.Verify(cache, time.Now()); err == nil {
		t.Fatal("the same announcement must not be accepted twice")
	}
}

func TestSignedAnnounceRejectsStaleTimestamp(t *testing.T) {
	id := testIdentity(t)
	sa, err := SignAnnounce(id, PeerInfo{Addr: "https://node-a.example:8080"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	future := time.Now().Add(announceMaxSkew + time.Minute)
	if err := sa.Verify(NewAnnounceNonceCache(), future); err == nil {
		t.Fatal("an announcement older than the skew window must be refused")
	}
}

func TestAnnounceNonceNotBurnedByBadSignature(t *testing.T) {
	id := testIdentity(t)
	cache := NewAnnounceNonceCache()
	sa, err := SignAnnounce(id, PeerInfo{Addr: "https://node-a.example:8080"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// An attacker who sees the nonce should not be able to spend it with a
	// forged signature and lock out the genuine announcement behind it.
	forged := *sa
	forged.Signature = append([]byte(nil), sa.Signature...)
	forged.Signature[0] ^= 0xff
	if err := forged.Verify(cache, time.Now()); err == nil {
		t.Fatal("forged signature should not verify")
	}

	if err := sa.Verify(cache, time.Now()); err != nil {
		t.Fatalf("genuine announcement must still be accepted: %v", err)
	}
}

func TestAnnounceNonceCacheExpires(t *testing.T) {
	cache := NewAnnounceNonceCache()
	nonce := make([]byte, announceNonceLen)
	nonce[0] = 1

	now := time.Now()
	if !cache.Use(nonce, now) {
		t.Fatal("first use should be accepted")
	}
	if cache.Use(nonce, now) {
		t.Fatal("immediate reuse should be rejected")
	}
	if !cache.Use(nonce, now.Add(announceNonceTTL+time.Second)) {
		t.Fatal("nonce should be forgotten after its TTL")
	}
}
