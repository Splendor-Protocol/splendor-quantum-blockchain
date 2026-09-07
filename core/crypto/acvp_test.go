package crypto

import (
	"bytes"
	"crypto/mlkem"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"testing"

	mldsa65 "github.com/cloudflare/circl/sign/mldsa/mldsa65"
)

// Conformance against the official NIST ACVP vectors for ML-DSA-65 (FIPS 204)
// and ML-KEM-768 (FIPS 203).
//
// The self-pinned vectors in mldsa_kat_test.go prove we did not CHANGE the
// scheme. These prove we implement the scheme NIST specified in the first
// place — a self-pinned vector would happily lock in a wrong answer forever.
//
// testdata/acvp-vectors.json is an unmodified subset of
// usnistgov/ACVP-Server, restricted to the two parameter sets this chain uses.

//go:embed testdata/acvp-vectors.json
var acvpVectorsJSON []byte

type acvpSuite struct {
	Source struct {
		Repo   string `json:"repo"`
		Commit string `json:"commit"`
	} `json:"_source"`

	MLDSAKeyGen []struct {
		TCID int    `json:"tcId"`
		Seed string `json:"seed"`
		PK   string `json:"pk"`
		SK   string `json:"sk"`
	} `json:"mldsa_keygen"`

	MLDSASigGen []struct {
		TCID      int    `json:"tcId"`
		SK        string `json:"sk"`
		Message   string `json:"message"`
		Context   string `json:"context"`
		Signature string `json:"signature"`
	} `json:"mldsa_siggen"`

	MLDSASigVer []struct {
		TCID       int    `json:"tcId"`
		PK         string `json:"pk"`
		Message    string `json:"message"`
		Context    string `json:"context"`
		Signature  string `json:"signature"`
		TestPassed bool   `json:"testPassed"`
		Reason     string `json:"reason"`
	} `json:"mldsa_sigver"`

	MLKEMKeyGen []struct {
		TCID int    `json:"tcId"`
		D    string `json:"d"`
		Z    string `json:"z"`
		EK   string `json:"ek"`
		DK   string `json:"dk"`
	} `json:"mlkem_keygen"`

	MLKEMEKCheck []struct {
		TCID       int    `json:"tcId"`
		EK         string `json:"ek"`
		TestPassed bool   `json:"testPassed"`
		Reason     string `json:"reason"`
	} `json:"mlkem_ek_check"`
}

func loadACVP(t *testing.T) *acvpSuite {
	t.Helper()
	var s acvpSuite
	if err := json.Unmarshal(acvpVectorsJSON, &s); err != nil {
		t.Fatalf("decode ACVP vectors: %v", err)
	}
	if len(s.MLDSAKeyGen) == 0 || len(s.MLDSASigGen) == 0 || len(s.MLKEMKeyGen) == 0 {
		t.Fatal("ACVP vector file is missing test groups")
	}
	return &s
}

func mustHex(t *testing.T, what, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decode %s: %v", what, err)
	}
	return b
}

// ── ML-DSA-65 (FIPS 204) ─────────────────────────────────────────────────────

// Key generation must reproduce NIST's keypair from NIST's seed.
func TestACVPMLDSAKeyGen(t *testing.T) {
	for _, v := range loadACVP(t).MLDSAKeyGen {
		seedBytes := mustHex(t, "seed", v.Seed)
		if len(seedBytes) != mldsa65.SeedSize {
			t.Fatalf("tc %d: seed is %d bytes, want %d", v.TCID, len(seedBytes), mldsa65.SeedSize)
		}
		var seed [mldsa65.SeedSize]byte
		copy(seed[:], seedBytes)

		pub, priv := mldsa65.NewKeyFromSeed(&seed)

		if !bytes.Equal((&PublicKey{k: pub}).Bytes(), mustHex(t, "pk", v.PK)) {
			t.Errorf("tc %d: public key does not match NIST", v.TCID)
		}
		if !bytes.Equal((&PrivateKey{k: priv}).Bytes(), mustHex(t, "sk", v.SK)) {
			t.Errorf("tc %d: private key does not match NIST", v.TCID)
		}
	}
}

// Deterministic signing must reproduce NIST's signature byte for byte.
//
// These vectors use the external, pure (non-pre-hashed) interface with a
// context string, which is precisely how this chain signs. Matching them is
// what makes the domain-separation contexts standards-conformant rather than
// merely self-consistent.
func TestACVPMLDSASigGen(t *testing.T) {
	for _, v := range loadACVP(t).MLDSASigGen {
		skBytes := mustHex(t, "sk", v.SK)
		msg := mustHex(t, "message", v.Message)
		ctx := mustHex(t, "context", v.Context)

		priv, err := PrivateKeyFromBytes(skBytes)
		if err != nil {
			t.Fatalf("tc %d: load private key: %v", v.TCID, err)
		}

		// Sign through the package API, so the wrapper is proven to be a
		// faithful pass-through and not quietly reshaping the input.
		sig, err := Sign(priv, msg, ctx)
		if err != nil {
			t.Fatalf("tc %d: sign: %v", v.TCID, err)
		}
		if !bytes.Equal(sig, mustHex(t, "signature", v.Signature)) {
			t.Errorf("tc %d: signature does not match NIST (context %d bytes, message %d bytes)",
				v.TCID, len(ctx), len(msg))
		}
	}
}

// Verification must agree with NIST on both the valid vector and each of the
// deliberately corrupted ones. Accepting a tampered signature here would mean
// accepting a forged transaction on-chain.
func TestACVPMLDSASigVer(t *testing.T) {
	for _, v := range loadACVP(t).MLDSASigVer {
		pub, err := PublicKeyFromBytes(mustHex(t, "pk", v.PK))
		if err != nil {
			t.Fatalf("tc %d: load public key: %v", v.TCID, err)
		}
		got := Verify(pub,
			mustHex(t, "message", v.Message),
			mustHex(t, "context", v.Context),
			mustHex(t, "signature", v.Signature),
		)
		if got != v.TestPassed {
			t.Errorf("tc %d (%s): Verify returned %v, NIST expects %v",
				v.TCID, v.Reason, got, v.TestPassed)
		}
	}
}

// ── ML-KEM-768 (FIPS 203) ────────────────────────────────────────────────────

// ML-KEM-768 key sizes, per FIPS 203, used to locate fields inside the
// expanded decapsulation key that NIST publishes.
const (
	mlkemDKPKESize768 = 1152 // dk_PKE
	mlkemHashSize     = 32
)

// Key generation must reproduce NIST's encapsulation key from NIST's (d, z).
//
// The standard library imports a decapsulation key only from its 64-byte seed
// and never exposes the 2400-byte expanded form, so NIST's dk cannot be loaded
// directly. Instead we check that the expanded key NIST published is consistent
// with the key we derived: FIPS 203 defines dk as dk_PKE || ek || H(ek) || z,
// so every field after dk_PKE is verifiable from our own output.
func TestACVPMLKEMKeyGen(t *testing.T) {
	for _, v := range loadACVP(t).MLKEMKeyGen {
		d := mustHex(t, "d", v.D)
		z := mustHex(t, "z", v.Z)
		dkExpanded := mustHex(t, "dk", v.DK)

		seed := append(append([]byte{}, d...), z...)
		if len(seed) != mlkem.SeedSize {
			t.Fatalf("tc %d: d||z is %d bytes, want %d", v.TCID, len(seed), mlkem.SeedSize)
		}

		dk, err := mlkem.NewDecapsulationKey768(seed)
		if err != nil {
			t.Fatalf("tc %d: key from seed: %v", v.TCID, err)
		}
		ek := dk.EncapsulationKey().Bytes()

		if !bytes.Equal(ek, mustHex(t, "ek", v.EK)) {
			t.Errorf("tc %d: encapsulation key does not match NIST", v.TCID)
			continue
		}

		if want := len(dkExpanded); want != mlkemDKPKESize768+len(ek)+mlkemHashSize+len(z) {
			t.Fatalf("tc %d: NIST dk is %d bytes, which does not match the FIPS 203 layout", v.TCID, want)
		}
		embeddedEK := dkExpanded[mlkemDKPKESize768 : mlkemDKPKESize768+len(ek)]
		if !bytes.Equal(embeddedEK, ek) {
			t.Errorf("tc %d: our encapsulation key is not the one embedded in NIST's dk", v.TCID)
		}
		hashOff := mlkemDKPKESize768 + len(ek)
		wantHash := Hash32(ek)
		if !bytes.Equal(dkExpanded[hashOff:hashOff+mlkemHashSize], wantHash[:]) {
			t.Errorf("tc %d: H(ek) inside NIST's dk does not match SHA3-256 of our ek", v.TCID)
		}
		if !bytes.Equal(dkExpanded[hashOff+mlkemHashSize:], z) {
			t.Errorf("tc %d: implicit-rejection value z inside NIST's dk does not match the input", v.TCID)
		}

		// An official key must also work end to end with our own code path.
		shared, ct, err := encapsulate(ek)
		if err != nil {
			t.Fatalf("tc %d: encapsulate to NIST key: %v", v.TCID, err)
		}
		recovered, err := dk.Decapsulate(ct)
		if err != nil {
			t.Fatalf("tc %d: decapsulate: %v", v.TCID, err)
		}
		if !bytes.Equal(shared, recovered) {
			t.Errorf("tc %d: shared secrets disagree for a NIST-derived key", v.TCID)
		}
	}
}

// Malformed encapsulation keys must be rejected. FIPS 203 requires the
// modulus check on import; skipping it would let a peer hand us a key that
// leaks information about the shared secret.
func TestACVPMLKEMEncapsulationKeyCheck(t *testing.T) {
	for _, v := range loadACVP(t).MLKEMEKCheck {
		_, err := mlkem.NewEncapsulationKey768(mustHex(t, "ek", v.EK))
		accepted := err == nil
		if accepted != v.TestPassed {
			t.Errorf("tc %d (%s): key accepted = %v, NIST expects %v",
				v.TCID, v.Reason, accepted, v.TestPassed)
		}
	}
}

// The vector file must stay traceable to its upstream source, so a future
// reader can re-derive it rather than trusting a blob in the repo.
func TestACVPVectorProvenance(t *testing.T) {
	s := loadACVP(t)
	if s.Source.Repo == "" || s.Source.Commit == "" {
		t.Fatal("ACVP vector file has no recorded upstream repo and commit")
	}
}
