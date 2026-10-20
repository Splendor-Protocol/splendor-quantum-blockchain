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

// Version 1 left from/to/amount/token outside the digest, so a signed
// settlement could be re-pointed at any recipient or amount. Version 2 binds
// all of them: changing any one after signing must fail both signatures, and
// a version-1 instruction is refused outright.
func TestVerifyInstructionBindsEverySettlementField(t *testing.T) {
	_, mldsaPriv, err := qcrypto.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_, slhPriv, err := qcrypto.GenerateSLHKeypair()
	if err != nil {
		t.Fatal(err)
	}
	ins := sampleInstruction()
	ins.SettlementAction = "native_transfer"
	ins.From = "SQ:" + repeatHex("11", 32)
	ins.To = "SQ:" + repeatHex("22", 32)
	ins.Amount = "1000000000000000000"
	ins.Token = "SQ:" + repeatHex("33", 32)
	if err := SignVerifyInstruction(DefaultChainID, ins, mldsaPriv, slhPriv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, _, err := CheckVerifyInstruction(DefaultChainID, ins); err != nil {
		t.Fatalf("check: %v", err)
	}

	tamper := map[string]func(*VerifyInstruction){
		"from":   func(x *VerifyInstruction) { x.From = "SQ:" + repeatHex("aa", 32) },
		"to":     func(x *VerifyInstruction) { x.To = "SQ:" + repeatHex("bb", 32) },
		"amount": func(x *VerifyInstruction) { x.Amount = "1000000000000000001" },
		"token":  func(x *VerifyInstruction) { x.Token = "SQ:" + repeatHex("cc", 32) },
		"action": func(x *VerifyInstruction) { x.SettlementAction = "qrc20_transfer" },
		"chain":  nil, // checked against another chain id below
	}
	for field, mut := range tamper {
		x := *ins
		chain := DefaultChainID
		if mut != nil {
			mut(&x)
		} else {
			chain = "splendor-quantum-testnet-1"
		}
		if _, _, err := CheckVerifyInstruction(chain, &x); err == nil {
			t.Fatalf("instruction with a tampered %s still verified", field)
		}
	}

	// Moving bytes between two adjacent fields must not collide: "ab"+"c"
	// and "a"+"bc" are different instructions.
	p := sampleInstruction()
	p.From, p.To = "ab", "c"
	q := sampleInstruction()
	q.From, q.To = "a", "bc"
	hp, _ := VerifyInstructionDigest(DefaultChainID, p)
	hq, _ := VerifyInstructionDigest(DefaultChainID, q)
	if hp == hq {
		t.Fatal("field boundaries are not bound: from/to bytes shifted without changing the digest")
	}

	// Version 1 is refused.
	v1 := sampleInstruction()
	v1.Version = 1
	if _, err := VerifyInstructionDigest(DefaultChainID, v1); err == nil {
		t.Fatal("a version-1 instruction was accepted")
	}
	// The digest prefix names the version.
	if string(verifyDigestPrefix) != "sqvl:instruction:v2:" {
		t.Fatalf("digest prefix = %q", verifyDigestPrefix)
	}
}

func repeatHex(pair string, n int) string {
	out := make([]byte, 0, 2*n)
	for i := 0; i < n; i++ {
		out = append(out, pair...)
	}
	return string(out)
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
