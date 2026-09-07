package types

import (
	"testing"

	qcrypto "github.com/splendor-protocol/splendor-quantum-blockchain/core/crypto"
)

func sampleInstruction() *VerifyInstruction {
	var id [32]byte
	id[0] = 0x11
	return &VerifyInstruction{
		Version:          VerifyInstructionVersion,
		InstructionID:    id,
		Purpose:          "payment",
		Origin:           "bank:test",
		Payload:          []byte("hello"),
		SettlementHint:   "splendor-qc",
		SettlementAction: "finalize_only",
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
	if _, _, err := CheckVerifyInstruction(DefaultChainID, ins); err != nil {
		t.Fatalf("check: %v", err)
	}

	// ML-DSA-only must fail (strip SLH).
	broken := *ins
	broken.SLHSig = broken.SLHSig[:len(broken.SLHSig)-1]
	if _, _, err := CheckVerifyInstruction(DefaultChainID, &broken); err == nil {
		t.Fatal("accepted an instruction with a truncated SLH-DSA signature")
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
