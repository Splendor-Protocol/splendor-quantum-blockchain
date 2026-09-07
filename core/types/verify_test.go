package types

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

func sampleInstruction() *VerifyInstruction {
	var id InstructionID
	id[0] = 0x11
	return &VerifyInstruction{
		Version:          VerifyInstructionVersion,
		InstructionID:    id,
		Purpose:          "payment",
		Origin:           "bank:test",
		Payload:          []byte("hello"),
		SettlementHint:   "splendor-qc",
		SettlementAction: SettleFinalizeOnly,
	}
}

func TestVerifyInstructionDualSigAndDomainSeparation(t *testing.T) {
	_, mldsaPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_, slhPriv, err := qcrypto.GenerateSLHKeypair()
	if err != nil {
		t.Fatal(err)
	}
	ins := sampleInstruction()
	if err := SignVerifyInstruction(DefaultChainID, ins, mldsaPriv, slhPriv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	_, signer, err := CheckVerifyInstruction(DefaultChainID, ins)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if signer != qcrypto.DeriveAddress(mldsaPriv.Public()) {
		t.Fatal("signer is not the ML-DSA key's address")
	}

	// ML-DSA-only must fail (strip SLH).
	broken := *ins
	broken.SLHSig = broken.SLHSig[:len(broken.SLHSig)-1]
	if _, _, err := CheckVerifyInstruction(DefaultChainID, &broken); err == nil {
		t.Fatal("accepted an instruction with a truncated SLH-DSA signature")
	}

	// Signed for one chain, presented on another.
	if _, _, err := CheckVerifyInstruction("splendor-quantum-other", ins); err == nil {
		t.Fatal("an instruction signed for one chain verified on another")
	}

	digest, err := VerifyInstructionDigest(DefaultChainID, ins)
	if err != nil {
		t.Fatal(err)
	}
	pk := mldsaPriv.Public()
	if qcrypto.Verify(pk, digest[:], qcrypto.CtxTransaction, ins.MLDSASig) {
		t.Fatal("instruction ML-DSA verified as a transaction signature")
	}
}

func TestVerifyInstructionDigestStable(t *testing.T) {
	a := sampleInstruction()
	b := sampleInstruction()
	ha, err := VerifyInstructionDigest(DefaultChainID, a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := VerifyInstructionDigest(DefaultChainID, b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatal("identical instructions produced different digests")
	}
	b.Origin = "bitcoin"
	hc, err := VerifyInstructionDigest(DefaultChainID, b)
	if err != nil {
		t.Fatal(err)
	}
	if ha == hc {
		t.Fatal("origin change did not change digest")
	}
}

// The id is hex on the wire in both directions, and the accidental
// encoding (an array of 32 integers) is refused rather than tolerated.
func TestInstructionIDIsHexOnTheWire(t *testing.T) {
	ins := sampleInstruction()
	raw, err := json.Marshal(ins)
	if err != nil {
		t.Fatal(err)
	}
	want := `"instruction_id":"1100000000000000000000000000000000000000000000000000000000000000"`
	if !bytes.Contains(raw, []byte(want)) {
		t.Fatalf("marshalled instruction lacks the hex id:\n%s", raw)
	}

	var back VerifyInstruction
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.InstructionID != ins.InstructionID {
		t.Fatal("hex id did not round-trip")
	}

	prefixed := strings.Replace(string(raw), `"instruction_id":"11`, `"instruction_id":"0x11`, 1)
	if err := json.Unmarshal([]byte(prefixed), &back); err != nil {
		t.Fatalf("0x-prefixed id refused: %v", err)
	}

	for name, in := range map[string]string{
		"array":    `{"instruction_id":[17,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]}`,
		"short":    `{"instruction_id":"1100"}`,
		"not hex":  `{"instruction_id":"zz00000000000000000000000000000000000000000000000000000000000000"}`,
		"a number": `{"instruction_id":17}`,
	} {
		var v VerifyInstruction
		if err := json.Unmarshal([]byte(in), &v); err == nil {
			t.Errorf("%s: an invalid instruction_id was accepted", name)
		}
	}

	if _, err := ParseInstructionID(strings.Repeat("ab", 32)); err != nil {
		t.Fatalf("64 hex chars refused: %v", err)
	}
	if got := ins.InstructionID.String(); len(got) != 64 {
		t.Fatalf("String() = %q", got)
	}
}

// An attestation is bound to the instance it was given to.
func TestVerifyAttestationDigestBindsTheInstance(t *testing.T) {
	var digest [32]byte
	digest[3] = 7
	a := qcrypto.Address{0xA}
	b := qcrypto.Address{0xB}
	da, err := VerifyAttestationDigest(DefaultChainID, a, digest)
	if err != nil {
		t.Fatal(err)
	}
	db, err := VerifyAttestationDigest(DefaultChainID, b, digest)
	if err != nil {
		t.Fatal(err)
	}
	if da == db {
		t.Fatal("attestation digest does not depend on the instance")
	}
	if _, err := VerifyAttestationDigest(DefaultChainID, qcrypto.Address{}, digest); err == nil {
		t.Fatal("zero instance accepted")
	}

	_, mldsaPriv, _ := qcrypto.GenerateKeypair()
	_, slhPriv, _ := qcrypto.GenerateSLHKeypair()
	sigM, sigS, err := SignVerifyAttestation(DefaultChainID, a, digest, mldsaPriv, slhPriv)
	if err != nil {
		t.Fatal(err)
	}
	if !qcrypto.Verify(mldsaPriv.Public(), da[:], qcrypto.CtxVerify, sigM) {
		t.Fatal("ML-DSA attestation signature does not verify over the attestation digest")
	}
	if !qcrypto.VerifySLH(slhPriv.Public(), da[:], qcrypto.CtxVerify, sigS) {
		t.Fatal("SLH-DSA attestation signature does not verify over the attestation digest")
	}
	if qcrypto.Verify(mldsaPriv.Public(), db[:], qcrypto.CtxVerify, sigM) {
		t.Fatal("attestation for instance A verified for instance B")
	}
}
